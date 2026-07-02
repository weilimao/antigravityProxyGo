import os
from bs4 import BeautifulSoup

def main():
    html_file = 'index.old.html'
    if not os.path.exists(html_file):
        print(f"File {html_file} not found!")
        return

    with open(html_file, 'r', encoding='utf-8') as f:
        content = f.read()

    soup = BeautifulSoup(content, 'html.parser')

    views = {
        'Dashboard': 'view-dashboard',
        'Accounts': 'view-accounts',
        'OTP': 'view-otp',
        'Settings': 'view-settings',
        'Packets': 'view-packets'
    }

    # Extract all top level modals to include them in the relevant views
    # For now, let's just create the basic Vue components.
    
    os.makedirs('src/views', exist_ok=True)
    
    for name, view_id in views.items():
        div = soup.find('div', id=view_id)
        if div:
            # Clean up class "hidden" so it displays correctly in Vue Router
            classes = div.get('class', [])
            if 'hidden' in classes:
                classes.remove('hidden')
            div['class'] = classes
            
            html_content = str(div)
            vue_content = f"""<template>
{html_content}
</template>

<script setup lang="ts">
import {{ onMounted }} from 'vue';

onMounted(() => {{
  // Logic from {name.lower()} controller will go here
}});
</script>
"""
            with open(f'src/views/{name}.vue', 'w', encoding='utf-8') as f:
                f.write(vue_content)
            print(f"Created {name}.vue")
        else:
            print(f"View {view_id} not found!")

if __name__ == '__main__':
    main()
